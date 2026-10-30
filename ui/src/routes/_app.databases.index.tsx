import { createFileRoute, redirect } from '@tanstack/react-router'

// The old address keeps working and keeps its filters.
export const Route=createFileRoute('/_app/databases/')({beforeLoad:({search}) => {throw redirect({to:'/data',search:(search) as never,replace:true})}})
