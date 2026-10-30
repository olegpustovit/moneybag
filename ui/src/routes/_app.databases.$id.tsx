import { createFileRoute, redirect } from '@tanstack/react-router'

export const Route=createFileRoute('/_app/databases/$id')({beforeLoad:({params}) => {throw redirect({to:'/data/$id',params,replace:true})}})
