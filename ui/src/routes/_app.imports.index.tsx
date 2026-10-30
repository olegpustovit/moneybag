import { createFileRoute, redirect } from '@tanstack/react-router'

// The old address keeps working and keeps its filters.
export const Route=createFileRoute('/_app/imports/')({beforeLoad:({search}) => {throw redirect({to:'/data/imports',search:(search) as never,replace:true})}})
