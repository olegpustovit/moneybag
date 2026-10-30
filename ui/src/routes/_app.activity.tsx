import { createFileRoute, redirect } from '@tanstack/react-router'

// The old address keeps working and keeps its filters.
export const Route=createFileRoute('/_app/activity')({beforeLoad:({search}) => {throw redirect({to:'/changes',search:(search) as never,replace:true})}})
